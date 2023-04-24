#!/home/edd1e/scripts/projs/uts_bot/uts_bot_env/bin/python3

import configuration as conf
from time import sleep
import os
import logging

from selenium import webdriver
from selenium.webdriver.chrome.service import Service
from selenium.webdriver.common.by import By
from selenium.webdriver.common.keys import Keys
from selenium.webdriver.support.ui import WebDriverWait
from selenium.webdriver.support import expected_conditions as EC
from selenium.common.exceptions import NoSuchElementException, StaleElementReferenceException,WebDriverException
from dotenv import load_dotenv
load_dotenv()

logging.basicConfig(
    level=logging.INFO, 
    format='%(asctime)s [%(levelname)s|%(name)s|%(funcName)s]:: %(message)s', 
    handlers=[logging.StreamHandler()]
)
class Browser:

    browser, service = None, None
    def __init__(self, driver:str):
        logging.info("INITIALIZING")
        web_options = webdriver.ChromeOptions() 
        web_options.add_argument("--remote-debugging-port=9222")  # thisself.service = Service(driver)
        web_options.add_experimental_option("detach", True)
        self.browser = webdriver.Chrome(service=self.service,options=web_options)
        self.browser.set_page_load_timeout(30)
        self.wait = WebDriverWait(self.browser, 30)
        

    def open_page(self,url:str):
        logging.info(f"OPENING:: {url}")
        self.browser.get(url)

    def click_button(self, element:object):
        try:
            element_to_string = Browser.get_attribute(element)
            logging.info(f"CLICKING ON ELEMENT:: {element_to_string}")
            element.click()
            return True
        except StaleElementReferenceException:
            logging.warning(f"StaleElementReferenceException, ELEMENT {element}, was not found")
            return False

    def find_inner_element(self,by_param:str, element:str, is_single_element=True):
        # if element == "section course-section":
        #     return self.browser.find_element(by_param, element)

        self.wait.until(EC.presence_of_element_located((by_param, element)))
        if is_single_element:
            return self.browser.find_element(by_param, element)
        else:
            return self.browser.find_elements(by_param, element)

    @classmethod
    def get_attribute(cls, element:object):
        try:
            str_element = str(element.get_attribute("class"))
            return str_element
        except StaleElementReferenceException:
            return "UKNOWN"

    def type_data(self, email_form:str, passwd_form:str, email:str, passwd:str):
        try:
            logging.info(f"TYPING DATA ON:: {email_form, passwd_form}")
            self.wait.until(EC.presence_of_element_located((By.NAME, email_form)))
            self.browser.find_element(By.NAME, email_form).send_keys(email + Keys.ENTER)
            sleep(1)
            self.wait.until(EC.presence_of_element_located((By.NAME, passwd_form)))
            self.browser.find_element(By.NAME, passwd_form).send_keys(passwd + Keys.ENTER)
        except NoSuchElementException:
            logging.warning("NoSuchElementException, trying again...")
            self.type_data(email_form, passwd_form, email, passwd)

    def go_back(self):
        # Navigate back and handle errors
        previous_url = self.browser.current_url
        try:
            logging.info("GOING TO PREVIOUS PAGE")
            self.browser.back()
        except WebDriverException as webExp:
            logging.warning("ERROR:: ", webExp)
            sleep(5) # Wait for some time
            if self.browser.current_url == previous_url:
                self.browser.refresh() # Refresh the page
                self.browser.back() # Try navigating back again
            logging.warning("TRYING AGAIN... GOING TO PREVIOUS PAGE")
            self.browser.back()


    def close_browser(self):
        logging.info("CLOSING BROWSER")
        self.browser.close()



if __name__ == "__main__":

    browser = Browser(conf.CHROME_DRIVER_DIR)
    browser.open_page(conf.saia_page)
    browser.click_button(browser.find_inner_element(By.CLASS_NAME, "login-identityprovider-btn"))
    browser.type_data("loginfmt", "passwd")
    browser.click_button(browser.find_inner_element(By.ID,"idBtn_Back"))
    browser.click_button(browser.find_inner_element(By.CLASS_NAME,'primary-navigation').find_elements(By.TAG_NAME, "li")[2].find_element(By.TAG_NAME, 'a'))
    sleep(2)
    courses = browser.find_inner_element(By.CLASS_NAME,"dashboard-card-deck").find_elements(By.CLASS_NAME,"dashboard-card")

    for element in courses:
        browser.click_button(element.find_element(By.TAG_NAME, "a"))
        sleep(2)
        browser.go_back()
    
    # browser.click_button(By.LINK_TEXT, "https://saia2.uts.edu.ve/my/courses.php")
    # browser.close_browser()